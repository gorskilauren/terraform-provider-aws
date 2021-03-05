package aws

import (
	"bytes"
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/mq"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/mitchellh/copystructure"
	"github.com/terraform-providers/terraform-provider-aws/aws/internal/hashcode"
	"github.com/terraform-providers/terraform-provider-aws/aws/internal/keyvaluetags"
)

func resourceAwsMqBroker() *schema.Resource {
	return &schema.Resource{
		Create: resourceAwsMqBrokerCreate,
		Read:   resourceAwsMqBrokerRead,
		Update: resourceAwsMqBrokerUpdate,
		Delete: resourceAwsMqBrokerDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		Schema: map[string]*schema.Schema{
			"apply_immediately": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"arn": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"authentication_strategy": {
				Type:         schema.TypeString,
				Optional:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringInSlice(mq.AuthenticationStrategy_Values(), true),
			},
			"auto_minor_version_upgrade": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
				ForceNew: true,
			},
			"broker_name": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"configuration": {
				Type:     schema.TypeList,
				Optional: true,
				Computed: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"id": {
							Type:     schema.TypeString,
							Optional: true,
							Computed: true,
						},
						"revision": {
							Type:     schema.TypeInt,
							Optional: true,
							Computed: true,
						},
					},
				},
			},
			"deployment_mode": {
				Type:         schema.TypeString,
				Optional:     true,
				Default:      mq.DeploymentModeSingleInstance,
				ForceNew:     true,
				ValidateFunc: validation.StringInSlice(mq.DeploymentMode_Values(), true),
			},
			"encryption_options": {
				Type:             schema.TypeList,
				Optional:         true,
				ForceNew:         true,
				MaxItems:         1,
				DiffSuppressFunc: suppressMissingOptionalConfigurationBlock,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"kms_key_id": {
							Type:         schema.TypeString,
							Optional:     true,
							Computed:     true,
							ForceNew:     true,
							ValidateFunc: validateArn,
						},
						"use_aws_owned_key": {
							Type:     schema.TypeBool,
							Optional: true,
							ForceNew: true,
							Default:  true,
						},
					},
				},
			},
			"engine_type": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringInSlice(mq.EngineType_Values(), true),
			},
			"engine_version": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"host_instance_type": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"instances": {
				Type:     schema.TypeList,
				Computed: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"console_url": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"endpoints": {
							Type:     schema.TypeList,
							Computed: true,
							Elem:     &schema.Schema{Type: schema.TypeString},
						},
						"ip_address": {
							Type:     schema.TypeString,
							Computed: true,
						},
					},
				},
			},
			"logs": {
				Type:     schema.TypeList,
				Optional: true,
				MaxItems: 1,
				// Ignore missing configuration block
				DiffSuppressFunc: func(k, old, new string, d *schema.ResourceData) bool {
					if old == "1" && new == "0" {
						return true
					}
					return false
				},
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"general": {
							Type:     schema.TypeBool,
							Optional: true,
						},
						"audit": {
							Type:     schema.TypeBool,
							Optional: true,
						},
					},
				},
			},
			"maintenance_window_start_time": {
				Type:     schema.TypeList,
				MaxItems: 1,
				Optional: true,
				Computed: true,
				ForceNew: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"day_of_week": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validation.StringInSlice(mq.DayOfWeek_Values(), true),
						},
						"time_of_day": {
							Type:     schema.TypeString,
							Required: true,
						},
						"time_zone": {
							Type:     schema.TypeString,
							Required: true,
						},
					},
				},
			},
			"publicly_accessible": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
				ForceNew: true,
			},
			"security_groups": {
				Type:     schema.TypeSet,
				Elem:     &schema.Schema{Type: schema.TypeString},
				Optional: true,
			},
			"storage_type": {
				Type:         schema.TypeString,
				Optional:     true,
				ValidateFunc: validation.StringInSlice(mq.BrokerStorageType_Values(), false),
			},
			"subnet_ids": {
				Type:     schema.TypeSet,
				Elem:     &schema.Schema{Type: schema.TypeString},
				Optional: true,
				Computed: true,
				ForceNew: true,
			},
			"tags": tagsSchema(),
			"user": {
				Type:     schema.TypeSet,
				Required: true,
				Set:      resourceAwsMqUserHash,
				DiffSuppressFunc: func(k, old, new string, d *schema.ResourceData) bool {
					// AWS currently does not support updating the RabbitMQ users beyond resource creation.
					// User list is not returned back after creation.
					// Updates to users can only be in the RabbitMQ UI.
					if v := d.Get("engine_type").(string); strings.EqualFold(v, mq.EngineTypeRabbitmq) && d.Get("arn").(string) != "" {
						return true
					}

					return false
				},
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"console_access": {
							Type:     schema.TypeBool,
							Optional: true,
							Default:  false,
						},
						"groups": {
							Type:     schema.TypeSet,
							MaxItems: 20,
							Elem: &schema.Schema{
								Type:         schema.TypeString,
								ValidateFunc: validation.StringLenBetween(2, 100),
							},
							Set:      schema.HashString,
							Optional: true,
						},
						"password": {
							Type:         schema.TypeString,
							Required:     true,
							Sensitive:    true,
							ValidateFunc: validateMqBrokerPassword,
						},
						"username": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validation.StringLenBetween(2, 100),
						},
					},
				},
			},
		},
	}
}

func resourceAwsMqBrokerCreate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).mqconn

	name := d.Get("broker_name").(string)
	requestId := resource.PrefixedUniqueId(fmt.Sprintf("tf-%s", name))
	input := mq.CreateBrokerRequest{
		AutoMinorVersionUpgrade: aws.Bool(d.Get("auto_minor_version_upgrade").(bool)),
		BrokerName:              aws.String(name),
		CreatorRequestId:        aws.String(requestId),
		EncryptionOptions:       expandMqEncryptionOptions(d.Get("encryption_options").([]interface{})),
		EngineType:              aws.String(d.Get("engine_type").(string)),
		EngineVersion:           aws.String(d.Get("engine_version").(string)),
		HostInstanceType:        aws.String(d.Get("host_instance_type").(string)),
		Logs:                    expandMqLogs(d.Get("logs").([]interface{})),
		PubliclyAccessible:      aws.Bool(d.Get("publicly_accessible").(bool)),
		Users:                   expandMqUsers(d.Get("user").(*schema.Set).List()),
	}

	if v, ok := d.GetOk("authentication_strategy"); ok {
		input.AuthenticationStrategy = aws.String(v.(string))
	}
	if v, ok := d.GetOk("configuration"); ok {
		input.Configuration = expandMqConfigurationId(v.([]interface{}))
	}
	if v, ok := d.GetOk("deployment_mode"); ok {
		input.DeploymentMode = aws.String(v.(string))
	}
	if v, ok := d.GetOk("maintenance_window_start_time"); ok {
		input.MaintenanceWindowStartTime = expandMqWeeklyStartTime(v.([]interface{}))
	}
	if v, ok := d.GetOk("security_groups"); ok && v.(*schema.Set).Len() > 0 {
		input.SecurityGroups = expandStringSet(d.Get("security_groups").(*schema.Set))
	}
	if v, ok := d.GetOk("storage_type"); ok {
		input.StorageType = aws.String(v.(string))
	}
	if v, ok := d.GetOk("subnet_ids"); ok {
		input.SubnetIds = expandStringSet(v.(*schema.Set))
	}
	if v, ok := d.GetOk("tags"); ok {
		input.Tags = keyvaluetags.New(v.(map[string]interface{})).IgnoreAws().MqTags()
	}

	log.Printf("[INFO] Creating MQ Broker: %s", input)
	out, err := conn.CreateBroker(&input)
	if err != nil {
		return err
	}

	d.SetId(aws.StringValue(out.BrokerId))
	d.Set("arn", out.BrokerArn)

	stateConf := resource.StateChangeConf{
		Pending: []string{
			mq.BrokerStateCreationInProgress,
			mq.BrokerStateRebootInProgress,
		},
		Target:  []string{mq.BrokerStateRunning},
		Timeout: 30 * time.Minute,
		Refresh: func() (interface{}, string, error) {
			out, err := conn.DescribeBroker(&mq.DescribeBrokerInput{
				BrokerId: aws.String(d.Id()),
			})
			if err != nil {
				return 42, "", err
			}

			return out, *out.BrokerState, nil
		},
	}
	_, err = stateConf.WaitForState()
	if err != nil {
		return err
	}

	return resourceAwsMqBrokerRead(d, meta)
}

func resourceAwsMqBrokerRead(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).mqconn
	ignoreTagsConfig := meta.(*AWSClient).IgnoreTagsConfig

	log.Printf("[INFO] Reading MQ Broker: %s", d.Id())
	out, err := conn.DescribeBroker(&mq.DescribeBrokerInput{
		BrokerId: aws.String(d.Id()),
	})
	if err != nil {
		if isAWSErr(err, mq.ErrCodeNotFoundException, "") {
			log.Printf("[WARN] MQ Broker %q not found, removing from state", d.Id())
			d.SetId("")
			return nil
		}
		// API docs say a 404 can also return a 403
		if isAWSErr(err, mq.ErrCodeForbiddenException, "Forbidden") {
			log.Printf("[WARN] MQ Broker %q not found, removing from state", d.Id())
			d.SetId("")
			return nil
		}
		return err
	}

	d.Set("arn", out.BrokerArn)
	d.Set("authentication_strategy", out.AuthenticationStrategy)
	d.Set("auto_minor_version_upgrade", out.AutoMinorVersionUpgrade)
	d.Set("broker_name", out.BrokerName)
	d.Set("deployment_mode", out.DeploymentMode)
	d.Set("engine_type", out.EngineType)
	d.Set("engine_version", out.EngineVersion)
	d.Set("host_instance_type", out.HostInstanceType)
	d.Set("instances", flattenMqBrokerInstances(out.BrokerInstances))
	d.Set("publicly_accessible", out.PubliclyAccessible)
	d.Set("security_groups", aws.StringValueSlice(out.SecurityGroups))
	d.Set("storage_type", out.StorageType)
	d.Set("subnet_ids", aws.StringValueSlice(out.SubnetIds))

	if err := d.Set("configuration", flattenMqConfigurationId(out.Configurations.Current)); err != nil {
		return fmt.Errorf("error setting configuration: %w", err)
	}
	if err := d.Set("encryption_options", flattenMqEncryptionOptions(out.EncryptionOptions)); err != nil {
		return fmt.Errorf("error setting encryption_options: %w", err)
	}
	if err := d.Set("logs", flattenMqLogs(out.Logs)); err != nil {
		return fmt.Errorf("error setting logs: %w", err)
	}
	if err := d.Set("maintenance_window_start_time", flattenMqWeeklyStartTime(out.MaintenanceWindowStartTime)); err != nil {
		return fmt.Errorf("error setting maintenance_window_start_time: %w", err)
	}

	rawUsers := make([]*mq.User, len(out.Users))
	for i, u := range out.Users {
		uOut, err := conn.DescribeUser(&mq.DescribeUserInput{
			BrokerId: aws.String(d.Id()),
			Username: u.Username,
		})
		if err != nil {
			return err
		}

		rawUsers[i] = &mq.User{
			ConsoleAccess: uOut.ConsoleAccess,
			Groups:        uOut.Groups,
			Username:      uOut.Username,
		}
	}

	if err := d.Set("user", flattenMqUsers(rawUsers, d.Get("user").(*schema.Set).List())); err != nil {
		return fmt.Errorf("error setting user: %w", err)
	}
	if err := d.Set("tags", keyvaluetags.MqKeyValueTags(out.Tags).IgnoreAws().IgnoreConfig(ignoreTagsConfig).Map()); err != nil {
		return fmt.Errorf("error setting tags: %w", err)
	}

	return nil
}

func resourceAwsMqBrokerUpdate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).mqconn

	requiresReboot := false

	if d.HasChange("security_groups") {
		_, err := conn.UpdateBroker(&mq.UpdateBrokerRequest{
			BrokerId:       aws.String(d.Id()),
			SecurityGroups: expandStringSet(d.Get("security_groups").(*schema.Set)),
		})
		if err != nil {
			return fmt.Errorf("error updating MQ Broker (%s) security groups: %w", d.Id(), err)
		}
	}

	if d.HasChanges("configuration", "logs") {
		_, err := conn.UpdateBroker(&mq.UpdateBrokerRequest{
			BrokerId:      aws.String(d.Id()),
			Configuration: expandMqConfigurationId(d.Get("configuration").([]interface{})),
			Logs:          expandMqLogs(d.Get("logs").([]interface{})),
		})
		if err != nil {
			return fmt.Errorf("error updating MQ Broker (%s) configuration: %w", d.Id(), err)
		}
		requiresReboot = true
	}

	if d.HasChange("user") {
		o, n := d.GetChange("user")
		var err error
		// d.HasChange("user") always reports a change when running resourceAwsMqBrokerUpdate
		// updateAwsMqBrokerUsers needs to be called to know if changes to user are actually made
		var usersUpdated bool
		usersUpdated, err = updateAwsMqBrokerUsers(conn, d.Id(),
			o.(*schema.Set).List(), n.(*schema.Set).List())
		if err != nil {
			return fmt.Errorf("error updating MQ Broker (%s) user: %w", d.Id(), err)
		}

		if usersUpdated {
			requiresReboot = true
		}
	}

	if d.Get("apply_immediately").(bool) && requiresReboot {
		_, err := conn.RebootBroker(&mq.RebootBrokerInput{
			BrokerId: aws.String(d.Id()),
		})
		if err != nil {
			return fmt.Errorf("error rebooting MQ Broker (%s): %w", d.Id(), err)
		}

		stateConf := resource.StateChangeConf{
			Pending: []string{
				mq.BrokerStateRunning,
				mq.BrokerStateRebootInProgress,
			},
			Target:  []string{mq.BrokerStateRunning},
			Timeout: 30 * time.Minute,
			Refresh: func() (interface{}, string, error) {
				out, err := conn.DescribeBroker(&mq.DescribeBrokerInput{
					BrokerId: aws.String(d.Id()),
				})
				if err != nil {
					return 42, "", err
				}

				return out, *out.BrokerState, nil
			},
		}
		_, err = stateConf.WaitForState()
		if err != nil {
			return err
		}
	}

	if d.HasChange("tags") {
		o, n := d.GetChange("tags")

		if err := keyvaluetags.MqUpdateTags(conn, d.Get("arn").(string), o, n); err != nil {
			return fmt.Errorf("error updating MQ Broker (%s) tags: %w", d.Get("arn").(string), err)
		}
	}

	return nil
}

func resourceAwsMqBrokerDelete(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).mqconn

	log.Printf("[INFO] Deleting MQ Broker: %s", d.Id())
	_, err := conn.DeleteBroker(&mq.DeleteBrokerInput{
		BrokerId: aws.String(d.Id()),
	})
	if err != nil {
		return err
	}

	return waitForMqBrokerDeletion(conn, d.Id())
}

func resourceAwsMqUserHash(v interface{}) int {
	var buf bytes.Buffer

	m := v.(map[string]interface{})
	if ca, ok := m["console_access"]; ok {
		buf.WriteString(fmt.Sprintf("%t-", ca.(bool)))
	} else {
		buf.WriteString("false-")
	}
	if g, ok := m["groups"]; ok {
		buf.WriteString(fmt.Sprintf("%v-", g.(*schema.Set).List()))
	}
	if p, ok := m["password"]; ok {
		buf.WriteString(fmt.Sprintf("%s-", p.(string)))
	}
	buf.WriteString(fmt.Sprintf("%s-", m["username"].(string)))

	return hashcode.String(buf.String())
}

func waitForMqBrokerDeletion(conn *mq.MQ, id string) error {
	stateConf := resource.StateChangeConf{
		Pending: []string{
			mq.BrokerStateRunning,
			mq.BrokerStateRebootInProgress,
			mq.BrokerStateDeletionInProgress,
		},
		Target:  []string{""},
		Timeout: 30 * time.Minute,
		Refresh: func() (interface{}, string, error) {
			out, err := conn.DescribeBroker(&mq.DescribeBrokerInput{
				BrokerId: aws.String(id),
			})
			if err != nil {
				if isAWSErr(err, "NotFoundException", "") {
					return 42, "", nil
				}
				return 42, "", err
			}

			return out, *out.BrokerState, nil
		},
	}
	_, err := stateConf.WaitForState()
	return err
}

func updateAwsMqBrokerUsers(conn *mq.MQ, bId string, oldUsers, newUsers []interface{}) (bool, error) {
	// If there are any user creates/deletes/updates, updatedUsers will be set to true
	updatedUsers := false

	createL, deleteL, updateL, err := diffAwsMqBrokerUsers(bId, oldUsers, newUsers)
	if err != nil {
		return updatedUsers, err
	}

	for _, c := range createL {
		_, err := conn.CreateUser(c)
		updatedUsers = true
		if err != nil {
			return updatedUsers, err
		}
	}
	for _, d := range deleteL {
		_, err := conn.DeleteUser(d)
		updatedUsers = true
		if err != nil {
			return updatedUsers, err
		}
	}
	for _, u := range updateL {
		_, err := conn.UpdateUser(u)
		updatedUsers = true
		if err != nil {
			return updatedUsers, err
		}
	}

	return updatedUsers, nil
}

func diffAwsMqBrokerUsers(bId string, oldUsers, newUsers []interface{}) (
	cr []*mq.CreateUserRequest, di []*mq.DeleteUserInput, ur []*mq.UpdateUserRequest, e error) {

	existingUsers := make(map[string]interface{})
	for _, ou := range oldUsers {
		u := ou.(map[string]interface{})
		username := u["username"].(string)
		// Convert Set to slice to allow easier comparison
		if g, ok := u["groups"]; ok {
			groups := g.(*schema.Set).List()
			u["groups"] = groups
		}

		existingUsers[username] = u
	}

	for _, nu := range newUsers {
		// Still need access to the original map
		// because Set contents doesn't get copied
		// Likely related to https://github.com/mitchellh/copystructure/issues/17
		nuOriginal := nu.(map[string]interface{})

		// Create a mutable copy
		newUser, err := copystructure.Copy(nu)
		if err != nil {
			return cr, di, ur, err
		}

		newUserMap := newUser.(map[string]interface{})
		username := newUserMap["username"].(string)

		// Convert Set to slice to allow easier comparison
		var ng []interface{}
		if g, ok := nuOriginal["groups"]; ok {
			ng = g.(*schema.Set).List()
			newUserMap["groups"] = ng
		}

		if eu, ok := existingUsers[username]; ok {

			existingUserMap := eu.(map[string]interface{})

			if !reflect.DeepEqual(existingUserMap, newUserMap) {
				ur = append(ur, &mq.UpdateUserRequest{
					BrokerId:      aws.String(bId),
					ConsoleAccess: aws.Bool(newUserMap["console_access"].(bool)),
					Groups:        expandStringList(ng),
					Password:      aws.String(newUserMap["password"].(string)),
					Username:      aws.String(username),
				})
			}

			// Delete after processing, so we know what's left for deletion
			delete(existingUsers, username)
		} else {
			cur := &mq.CreateUserRequest{
				BrokerId:      aws.String(bId),
				ConsoleAccess: aws.Bool(newUserMap["console_access"].(bool)),
				Password:      aws.String(newUserMap["password"].(string)),
				Username:      aws.String(username),
			}
			if len(ng) > 0 {
				cur.Groups = expandStringList(ng)
			}
			cr = append(cr, cur)
		}
	}

	for username := range existingUsers {
		di = append(di, &mq.DeleteUserInput{
			BrokerId: aws.String(bId),
			Username: aws.String(username),
		})
	}

	return cr, di, ur, nil
}

func expandMqEncryptionOptions(l []interface{}) *mq.EncryptionOptions {
	if len(l) == 0 || l[0] == nil {
		return nil
	}

	m := l[0].(map[string]interface{})

	encryptionOptions := &mq.EncryptionOptions{
		UseAwsOwnedKey: aws.Bool(m["use_aws_owned_key"].(bool)),
	}

	if v, ok := m["kms_key_id"].(string); ok && v != "" {
		encryptionOptions.KmsKeyId = aws.String(v)
	}

	return encryptionOptions
}

func flattenMqEncryptionOptions(encryptionOptions *mq.EncryptionOptions) []interface{} {
	if encryptionOptions == nil {
		return []interface{}{}
	}

	m := map[string]interface{}{
		"kms_key_id":        aws.StringValue(encryptionOptions.KmsKeyId),
		"use_aws_owned_key": aws.BoolValue(encryptionOptions.UseAwsOwnedKey),
	}

	return []interface{}{m}
}

func validateMqBrokerPassword(v interface{}, k string) (ws []string, errors []error) {
	min := 12
	max := 250
	value := v.(string)
	unique := make(map[string]bool)

	for _, v := range value {
		if _, ok := unique[string(v)]; ok {
			continue
		}
		if string(v) == "," {
			errors = append(errors, fmt.Errorf("%q must not contain commas", k))
		}
		unique[string(v)] = true
	}
	if len(unique) < 4 {
		errors = append(errors, fmt.Errorf("%q must contain at least 4 unique characters", k))
	}
	if len(value) < min || len(value) > max {
		errors = append(errors, fmt.Errorf(
			"%q must be %d to %d characters long. provided string length: %d", k, min, max, len(value)))
	}
	return
}

func expandMqUsers(cfg []interface{}) []*mq.User {
	users := make([]*mq.User, len(cfg))
	for i, m := range cfg {
		u := m.(map[string]interface{})
		user := mq.User{
			Username: aws.String(u["username"].(string)),
			Password: aws.String(u["password"].(string)),
		}
		if v, ok := u["console_access"]; ok {
			user.ConsoleAccess = aws.Bool(v.(bool))
		}
		if v, ok := u["groups"]; ok {
			user.Groups = expandStringSet(v.(*schema.Set))
		}
		users[i] = &user
	}
	return users
}

// We use cfgdUsers to get & set the password
func flattenMqUsers(users []*mq.User, cfgUsers []interface{}) *schema.Set {
	existingPairs := make(map[string]string)
	for _, u := range cfgUsers {
		user := u.(map[string]interface{})
		username := user["username"].(string)
		existingPairs[username] = user["password"].(string)
	}

	out := make([]interface{}, 0)
	for _, u := range users {
		m := map[string]interface{}{
			"username": *u.Username,
		}
		password := ""
		if p, ok := existingPairs[*u.Username]; ok {
			password = p
		}
		if password != "" {
			m["password"] = password
		}
		if u.ConsoleAccess != nil {
			m["console_access"] = *u.ConsoleAccess
		}
		if len(u.Groups) > 0 {
			m["groups"] = flattenStringSet(u.Groups)
		}
		out = append(out, m)
	}
	return schema.NewSet(resourceAwsMqUserHash, out)
}

func expandMqWeeklyStartTime(cfg []interface{}) *mq.WeeklyStartTime {
	if len(cfg) < 1 {
		return nil
	}

	m := cfg[0].(map[string]interface{})
	return &mq.WeeklyStartTime{
		DayOfWeek: aws.String(m["day_of_week"].(string)),
		TimeOfDay: aws.String(m["time_of_day"].(string)),
		TimeZone:  aws.String(m["time_zone"].(string)),
	}
}

func flattenMqWeeklyStartTime(wst *mq.WeeklyStartTime) []interface{} {
	if wst == nil {
		return []interface{}{}
	}
	m := make(map[string]interface{})
	if wst.DayOfWeek != nil {
		m["day_of_week"] = *wst.DayOfWeek
	}
	if wst.TimeOfDay != nil {
		m["time_of_day"] = *wst.TimeOfDay
	}
	if wst.TimeZone != nil {
		m["time_zone"] = *wst.TimeZone
	}
	return []interface{}{m}
}

func expandMqConfigurationId(cfg []interface{}) *mq.ConfigurationId {
	if len(cfg) < 1 {
		return nil
	}

	m := cfg[0].(map[string]interface{})
	out := mq.ConfigurationId{
		Id: aws.String(m["id"].(string)),
	}
	if v, ok := m["revision"].(int); ok && v > 0 {
		out.Revision = aws.Int64(int64(v))
	}

	return &out
}

func flattenMqConfigurationId(cid *mq.ConfigurationId) []interface{} {
	if cid == nil {
		return []interface{}{}
	}
	m := make(map[string]interface{})
	if cid.Id != nil {
		m["id"] = *cid.Id
	}
	if cid.Revision != nil {
		m["revision"] = *cid.Revision
	}
	return []interface{}{m}
}

func flattenMqBrokerInstances(instances []*mq.BrokerInstance) []interface{} {
	if len(instances) == 0 {
		return []interface{}{}
	}
	l := make([]interface{}, len(instances))
	for i, instance := range instances {
		m := make(map[string]interface{})
		if instance.ConsoleURL != nil {
			m["console_url"] = *instance.ConsoleURL
		}
		if len(instance.Endpoints) > 0 {
			m["endpoints"] = aws.StringValueSlice(instance.Endpoints)
		}
		if instance.IpAddress != nil {
			m["ip_address"] = *instance.IpAddress
		}
		l[i] = m
	}

	return l
}

func flattenMqLogs(logs *mq.LogsSummary) []interface{} {
	if logs == nil {
		return []interface{}{}
	}

	m := map[string]interface{}{}

	if logs.General != nil {
		m["general"] = aws.BoolValue(logs.General)
	}

	if logs.Audit != nil {
		m["audit"] = aws.BoolValue(logs.Audit)
	}

	return []interface{}{m}
}

func expandMqLogs(l []interface{}) *mq.Logs {
	if len(l) == 0 || l[0] == nil {
		return nil
	}

	m := l[0].(map[string]interface{})

	logs := &mq.Logs{}

	if v, ok := m["general"]; ok {
		logs.General = aws.Bool(v.(bool))
	}

	if v, ok := m["audit"]; ok {
		logs.Audit = aws.Bool(v.(bool))
	}

	return logs
}
