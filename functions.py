#!/home/edd1e/scripts/projs/uts_bot/uts_bot_env/bin/python3
import configuration as conf
import driver
import helpers

from time import sleep
import os
import logging



from selenium.webdriver.common.by import By
from selenium.webdriver.common.keys import Keys
from dotenv import load_dotenv
load_dotenv()
logging.basicConfig(
    level=logging.INFO, 
    format='%(asctime)s [%(levelname)s|%(name)s|%(funcName)s]:: %(message)s', 
    handlers=[logging.StreamHandler()]
)
class Get_Saia_Data:


    def __init__(self, email, passwd):
        self.email = email
        self.passwd = passwd
        self.driver = driver.Browser(conf.CHROME_DRIVER_DIR)


    def get_courses_deadlines(self):
        #Enter saia page
        self.driver.open_page(conf.saia_page)

        #Enter login button
        self.driver.click_button(self.driver.find_inner_element(By.CLASS_NAME, "login-identityprovider-btn"))
        
        #Type in login and password data
        self.driver.type_data("loginfmt", "passwd", self.email, self.passwd)

        #Click on 'Not now' button after login
        self.driver.click_button(self.driver.find_inner_element(By.ID,"idBtn_Back"))

        #Click on 'Courses' button 
        self.driver.click_button(self.driver.find_inner_element(By.CLASS_NAME,'primary-navigation').find_elements(By.TAG_NAME, "li")[2].find_element(By.TAG_NAME, 'a'))
        
        courses = self.driver.find_inner_element(By.CLASS_NAME,"dashboard-card-deck").find_elements(By.CLASS_NAME,"dashboard-card")
        
        #Now for every course on dashboard, it will click it and enter it 
        for element in courses:
            get_activities_helper = helpers.Helpers(self.driver)
            get_activities_helper.get_saia_activities(element)
            self.driver.go_back()


if __name__ == "__main__":
    email = os.getenv('EMAIL')
    password = os.getenv('PASSWD')
    browser = Get_Saia_Data(email,password)
    browser.get_courses_deadlines()